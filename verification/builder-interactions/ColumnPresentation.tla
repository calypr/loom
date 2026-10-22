---- MODULE ColumnPresentation ----
EXTENDS Naturals, TLC

VARIABLES c, lastAction

vars == <<c, lastAction>>

TypeOK ==
  /\ c.label \in {"A", "B"}
  /\ c.visible \in BOOLEAN
  /\ c.order \in 0..2
  /\ c.filterOffered \in BOOLEAN
  /\ c.filterCapability \in BOOLEAN
  /\ c.featurePolicy \in {"NONE", "SUM", "MEAN", "FIRST_ORDERED"}
  /\ c.searchQuery \in {"", "a", "zz"}
  /\ c.searchResults \in 0..2
  /\ c.rowValuePolicy \in {"ONE", "ALL"}
  /\ c.inspectPhase \in {"idle", "loading", "ready", "rejected"}
  /\ c.inspectColumn \in {"NONE", "C1", "C2"}
  /\ c.inspectResponse \in {"NONE", "C1", "C2"}
  /\ c.relatedAggregatePhase \in {"idle", "created"}
  /\ c.removed \in BOOLEAN
  /\ lastAction \in {
       "Init", "RenameColumn", "MoveColumn", "ToggleVisibility",
       "SetFilterOffered", "SetFeaturePolicy", "SearchColumns",
       "SetRowValuePolicy", "InspectColumn", "InspectSucceeded",
       "InspectRejected", "CreateRelatedAggregate", "RemoveConfiguredColumn"
     }

Init ==
  /\ c = [
       label |-> "A",
       visible |-> TRUE,
       order |-> 0,
       filterOffered |-> FALSE,
       filterCapability |-> TRUE,
       featurePolicy |-> "NONE",
       searchQuery |-> "",
       searchResults |-> 2,
       rowValuePolicy |-> "ONE",
       inspectPhase |-> "idle",
       inspectColumn |-> "NONE",
       inspectResponse |-> "NONE",
       relatedAggregatePhase |-> "idle",
       removed |-> FALSE
     ]
  /\ lastAction = "Init"

RenameColumn(newLabel) ==
  /\ newLabel \in {"A", "B", ""}
  /\ c' = [c EXCEPT !.label = IF newLabel = "" THEN c.label ELSE newLabel]
  /\ lastAction' = "RenameColumn"

MoveColumn(newOrder) ==
  /\ newOrder \in 0..2
  /\ c' = [c EXCEPT !.order = newOrder]
  /\ lastAction' = "MoveColumn"

ToggleVisibility ==
  /\ ~c.removed
  /\ c' = [c EXCEPT !.visible = ~c.visible]
  /\ lastAction' = "ToggleVisibility"

SetFilterOffered(value) ==
  /\ value \in BOOLEAN
  /\ (value => c.filterCapability)
  /\ c' = [c EXCEPT !.filterOffered = value]
  /\ lastAction' = "SetFilterOffered"

SetFeaturePolicy(policy) ==
  /\ policy \in {"NONE", "SUM", "MEAN", "FIRST_ORDERED"}
  /\ c' = [c EXCEPT !.featurePolicy = policy]
  /\ lastAction' = "SetFeaturePolicy"

SearchColumns(query) ==
  /\ query \in {"", "a", "zz"}
  /\ c' = [c EXCEPT
       !.searchQuery = query,
       !.searchResults = IF query = "" THEN 2 ELSE IF query = "a" THEN 1 ELSE 0
     ]
  /\ lastAction' = "SearchColumns"

SetRowValuePolicy(policy) ==
  /\ policy \in {"ONE", "ALL"}
  /\ c' = [c EXCEPT !.rowValuePolicy = policy]
  /\ lastAction' = "SetRowValuePolicy"

InspectColumn(column) ==
  /\ column \in {"C1", "C2"}
  /\ c' = [c EXCEPT
       !.inspectPhase = "loading",
       !.inspectColumn = column,
       !.inspectResponse = "NONE"
     ]
  /\ lastAction' = "InspectColumn"

InspectSucceeded(response) ==
  /\ c.inspectPhase = "loading"
  /\ response = c.inspectColumn
  /\ c' = [c EXCEPT !.inspectPhase = "ready", !.inspectResponse = response]
  /\ lastAction' = "InspectSucceeded"

InspectRejected(response) ==
  /\ c.inspectPhase = "loading"
  /\ response # c.inspectColumn
  /\ c' = [c EXCEPT !.inspectPhase = "rejected", !.inspectResponse = response]
  /\ lastAction' = "InspectRejected"

CreateRelatedAggregate ==
  /\ c.relatedAggregatePhase = "idle"
  /\ c' = [c EXCEPT !.relatedAggregatePhase = "created"]
  /\ lastAction' = "CreateRelatedAggregate"

RemoveConfiguredColumn ==
  /\ c' = [c EXCEPT !.removed = TRUE, !.visible = FALSE]
  /\ lastAction' = "RemoveConfiguredColumn"

Next ==
  \/ \E newLabel \in {"A", "B", ""}: RenameColumn(newLabel)
  \/ \E newOrder \in 0..2: MoveColumn(newOrder)
  \/ ToggleVisibility
  \/ \E value \in BOOLEAN: SetFilterOffered(value)
  \/ \E policy \in {"NONE", "SUM", "MEAN", "FIRST_ORDERED"}: SetFeaturePolicy(policy)
  \/ \E query \in {"", "a", "zz"}: SearchColumns(query)
  \/ \E policy \in {"ONE", "ALL"}: SetRowValuePolicy(policy)
  \/ \E column \in {"C1", "C2"}: InspectColumn(column)
  \/ \E response \in {"C1", "C2"}: InspectSucceeded(response)
  \/ \E response \in {"NONE", "C1", "C2"}: InspectRejected(response)
  \/ CreateRelatedAggregate
  \/ RemoveConfiguredColumn

Spec == Init /\ [][Next]_vars

Inv_LabelNonEmpty ==
  c.label # ""

Inv_FilterRequiresCapability ==
  c.filterOffered => c.filterCapability

Inv_SearchResultsMatchQuery ==
  c.searchResults =
    (IF c.searchQuery = "" THEN 2 ELSE IF c.searchQuery = "a" THEN 1 ELSE 0)

Inv_ReadyInspectionMatchesColumn ==
  c.inspectPhase = "ready" =>
    /\ c.inspectResponse = c.inspectColumn
    /\ c.inspectColumn # "NONE"

Inv_RejectedInspectionMismatch ==
  c.inspectPhase = "rejected" => c.inspectResponse # c.inspectColumn

Inv_RemovedColumnsAreHidden ==
  c.removed => ~c.visible

Inv_PresentationBounds ==
  /\ c.order \in 0..2
  /\ c.rowValuePolicy \in {"ONE", "ALL"}
  /\ c.featurePolicy \in {"NONE", "SUM", "MEAN", "FIRST_ORDERED"}

====
