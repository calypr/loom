---------------- MODULE BuilderJourney ----------------
\* A composed, bounded user journey: select a row source, apply at most two
\* reshapes, optionally pivot, then preview the current table.
\*
\* The model deliberately keeps data shape out of scope. A trace observation
\* maps the rendered UI state onto phase/preview state. Data-shape correctness
\* remains in BuilderOperations.tla and the fixture oracle carried by the
\* browser trace.
EXTENDS Naturals, Sequences, TLC

MaxReshapes == 2

Phases == {"idle", "selected", "reshaping"}
PreviewStates == {"none", "pending", "ready", "error"}
JourneyActions == {"Init", "SelectRows", "Group", "GroupAgain", "AddColumn", "AddColumnAgain",
                   "Filter", "CancelFilter", "Unpivot", "EditGroup", "RemoveGroup", "Pivot", "PivotDirect", "Preview", "PreviewComplete",
                   "PreviewError"}

VARIABLES
  phase,
  reshapeCount,
  pivotReady,
  preview,
  rowsPresent,
  lastAction

vars == <<phase, reshapeCount, pivotReady, preview, rowsPresent, lastAction>>

Init ==
  /\ phase = "idle"
  /\ reshapeCount = 0
  /\ pivotReady = FALSE
  /\ preview = "none"
  /\ rowsPresent = FALSE
  /\ lastAction = "Init"

SelectRows ==
  /\ phase = "idle"
  /\ phase' = "selected"
  /\ UNCHANGED <<reshapeCount, pivotReady, preview, rowsPresent>>
  /\ lastAction' = "SelectRows"

Group ==
  /\ phase \in {"selected", "reshaping"}
  /\ ~pivotReady
  /\ reshapeCount < MaxReshapes
  /\ phase' = "reshaping"
  /\ reshapeCount' = reshapeCount + 1
  /\ UNCHANGED <<pivotReady, preview, rowsPresent>>
  /\ lastAction' = "Group"

GroupAgain ==
  /\ phase = "reshaping"
  /\ ~pivotReady
  /\ reshapeCount = 1
  /\ reshapeCount' = 2
  /\ UNCHANGED <<phase, pivotReady, preview, rowsPresent>>
  /\ lastAction' = "GroupAgain"

AddColumn ==
  /\ phase \in {"selected", "reshaping"}
  /\ ~pivotReady
  /\ reshapeCount < MaxReshapes
  /\ phase' = "reshaping"
  /\ reshapeCount' = reshapeCount + 1
  /\ UNCHANGED <<pivotReady, preview, rowsPresent>>
  /\ lastAction' = "AddColumn"

AddColumnAgain ==
  /\ phase = "reshaping"
  /\ ~pivotReady
  /\ reshapeCount = 1
  /\ reshapeCount' = 2
  /\ UNCHANGED <<phase, pivotReady, preview, rowsPresent>>
  /\ lastAction' = "AddColumnAgain"

Filter ==
  /\ phase \in {"selected", "reshaping"}
  /\ ~pivotReady
  /\ reshapeCount < MaxReshapes
  /\ phase' = "reshaping"
  /\ reshapeCount' = reshapeCount + 1
  /\ UNCHANGED <<pivotReady, preview, rowsPresent>>
  /\ lastAction' = "Filter"

CancelFilter ==
  /\ phase = "reshaping"
  /\ reshapeCount = 1
  /\ preview = "ready"
  /\ phase' = "selected"
  /\ reshapeCount' = 0
  /\ preview' = "none"
  /\ rowsPresent' = FALSE
  /\ UNCHANGED pivotReady
  /\ lastAction' = "CancelFilter"

Unpivot ==
  /\ phase \in {"selected", "reshaping"}
  /\ ~pivotReady
  /\ reshapeCount < MaxReshapes
  /\ phase' = "reshaping"
  /\ reshapeCount' = reshapeCount + 1
  /\ UNCHANGED <<pivotReady, preview, rowsPresent>>
  /\ lastAction' = "Unpivot"

EditGroup ==
  /\ phase = "reshaping"
  /\ reshapeCount = 1
  /\ ~pivotReady
  /\ reshapeCount' = 2
  /\ UNCHANGED <<phase, pivotReady, preview, rowsPresent>>
  /\ lastAction' = "EditGroup"

RemoveGroup ==
  /\ phase = "reshaping"
  /\ reshapeCount >= 1
  /\ ~pivotReady
  /\ phase' = "selected"
  /\ reshapeCount' = 0
  /\ preview' = "none"
  /\ rowsPresent' = FALSE
  /\ UNCHANGED pivotReady
  /\ lastAction' = "RemoveGroup"

Pivot ==
  /\ phase = "reshaping"
  /\ reshapeCount = MaxReshapes
  /\ ~pivotReady
  /\ pivotReady' = TRUE
  /\ UNCHANGED <<phase, reshapeCount, preview, rowsPresent>>
  /\ lastAction' = "Pivot"

PivotDirect ==
  /\ phase = "selected"
  /\ ~pivotReady
  /\ pivotReady' = TRUE
  /\ UNCHANGED <<phase, reshapeCount, preview, rowsPresent>>
  /\ lastAction' = "PivotDirect"

Preview ==
  /\ (pivotReady \/ reshapeCount > 0)
  /\ preview = "none"
  /\ preview' = "pending"
  /\ UNCHANGED <<phase, reshapeCount, pivotReady, rowsPresent>>
  /\ lastAction' = "Preview"

PreviewComplete ==
  /\ preview = "pending"
  /\ preview' = "ready"
  /\ rowsPresent' = TRUE
  /\ UNCHANGED <<phase, reshapeCount, pivotReady>>
  /\ lastAction' = "PreviewComplete"

PreviewError ==
  /\ preview = "pending"
  /\ preview' = "error"
  /\ rowsPresent' = FALSE
  /\ UNCHANGED <<phase, reshapeCount, pivotReady>>
  /\ lastAction' = "PreviewError"

Stutter ==
  UNCHANGED vars

Next ==
  \/ SelectRows
  \/ Group
  \/ GroupAgain
  \/ AddColumn
  \/ AddColumnAgain
  \/ Filter
  \/ CancelFilter
  \/ Unpivot
  \/ EditGroup
  \/ RemoveGroup
  \/ Pivot
  \/ PivotDirect
  \/ Preview
  \/ PreviewComplete
  \/ PreviewError
  \/ Stutter

Spec == Init /\ [][Next]_vars

TypeOK ==
  /\ phase \in Phases
  /\ reshapeCount \in 0..MaxReshapes
  /\ pivotReady \in BOOLEAN
  /\ preview \in PreviewStates
  /\ rowsPresent \in BOOLEAN
  /\ lastAction \in JourneyActions

\* A preview may follow at least one reshape or a direct pivot.
PreviewRequiresReshapeOrPivot ==
  preview \in {"pending", "ready", "error"} => (reshapeCount > 0 \/ pivotReady)

PivotRequiresReshapeOrDirect ==
  pivotReady => (reshapeCount = MaxReshapes \/ phase = "selected")

RowsOnlyAfterPreview ==
  rowsPresent => preview = "ready"

=============================================================================
