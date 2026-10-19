---------------- MODULE BuilderOperations ----------------
EXTENDS Naturals, FiniteSets, Sequences, TLC

(*
  Bounded operation/data-shape model for the mounted Builder editors.

  The model enumerates the construction operations the mounted UI offers and
  checks that every offered, well-formed operation is one the authoring
  backend accepts.  It also checks that every authored step receives exactly
  the columns emitted by its predecessor.

  This module deliberately contains no proposal/preview/apply concurrency.
  BuilderPreview.tla owns those asynchronous states.  Keeping the two finite
  models separate prevents an operation-sequence cross product from obscuring
  the lifecycle counterexamples.

  Source map:
    ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/
    ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx
    internal/explorer/authoringv2/construction_validation.go
    internal/explorer/lifecycle/construction_proposal.go

  Bound: two authored steps over the operation families the mounted editors
  expose for a rooted table.  TLC explores every reachable ordering in that
  bound.  This is not a proof over arbitrary construction depth.
*)

MaxSteps == 2
StepIds == {1, 2}
ColumnClasses == {"string", "integer", "list_string", "related_coding"}
ScalarClasses == {"string", "integer"}
NumericClasses == {"integer"}
BaseColumns == ColumnClasses
Shapes == {"records", "groups", "expanded"}
NoOp == "none"
NoStep == 0
LastValues == StepIds \union {NoStep}

Ops == {
  "ADD_FIELD", "ADD_RELATED", "FILTER", "RELATED_ELIGIBILITY", "DERIVE", "GROUP", "PIVOT",
  "CODED_PIVOT", "UNPIVOT", "EXPAND", "RELATED_EXPAND",
  "COMBINE_APPEND", "COMBINE_KEY_JOIN", "COMBINE_MEMBERSHIP",
  "ROW_EXPANDED", "ROW_EXPLICIT_GROUP",
  "TABLE_SHAPE_PIVOT", "TABLE_SHAPE_UNPIVOT"
}
OpValues == Ops \union {NoOp}
AuthorableOps == {
  "ADD_FIELD", "ADD_RELATED", "FILTER", "RELATED_ELIGIBILITY", "DERIVE", "GROUP", "PIVOT",
  "UNPIVOT", "EXPAND", "RELATED_EXPAND"
}
EditFamilies == {"NONE", "KEEP_ROWS", "CALCULATE", "RESHAPE", "COMBINE", "RELATED"}
EditFamily(op) ==
  CASE op = "FILTER" -> "KEEP_ROWS"
    [] op = "RELATED_ELIGIBILITY" -> "KEEP_ROWS"
    [] op = "DERIVE" -> "CALCULATE"
    [] op = "GROUP" -> "RESHAPE"
    [] op = "PIVOT" -> "RESHAPE"
    [] op = "CODED_PIVOT" -> "RESHAPE"
    [] op = "UNPIVOT" -> "RESHAPE"
    [] op = "EXPAND" -> "RESHAPE"
    [] op = "RELATED_EXPAND" -> "RELATED"
    [] op = "ADD_RELATED" -> "RELATED"
    [] op = "COMBINE_APPEND" -> "COMBINE"
    [] op = "COMBINE_KEY_JOIN" -> "COMBINE"
    [] op = "COMBINE_MEMBERSHIP" -> "COMBINE"
    [] OTHER -> "NONE"

ScalarCount(cols) == Cardinality(cols \intersect ScalarClasses)
NumericCount(cols) == Cardinality(cols \intersect NumericClasses)

(* Bounded backend support.  "unsupported" is a typed refusal of a valid form. *)
StepSupport(op, shape, cols, constructionPresent, stepCount, relatedAnchor) ==
  CASE op = "ADD_FIELD" -> "supported"
    [] op = "ADD_RELATED" ->
         IF "related_coding" \in cols THEN "supported" ELSE "unsupported"
    [] op = "FILTER" ->
         IF ScalarCount(cols) >= 1 THEN "supported" ELSE "unsupported"
    [] op = "RELATED_ELIGIBILITY" ->
         IF relatedAnchor /\ ScalarCount(cols) >= 1 THEN "supported" ELSE "unsupported"
    [] op = "DERIVE" ->
         IF NumericCount(cols) >= 1 THEN "supported" ELSE "unsupported"
    [] op = "GROUP" ->
         IF ScalarCount(cols) >= 1 THEN "supported" ELSE "unsupported"
    [] op = "PIVOT" ->
         IF ScalarCount(cols) >= 2 THEN "supported" ELSE "unsupported"
    [] op = "CODED_PIVOT" ->
         IF "code" \in cols THEN "supported" ELSE "unsupported"
    [] op = "UNPIVOT" ->
         IF ScalarCount(cols) >= 2 THEN "supported" ELSE "unsupported"
    [] op = "EXPAND" ->
         IF "list_string" \in cols /\ shape = "records" /\ stepCount < 2
         THEN "supported" ELSE "unsupported"
    [] op = "RELATED_EXPAND" ->
         IF "related_coding" \in cols THEN "supported" ELSE "unsupported"
    [] op = "COMBINE_APPEND" ->
         IF stepCount >= 1 THEN "supported" ELSE "unsupported"
    [] op = "COMBINE_KEY_JOIN" ->
         IF stepCount >= 1 THEN "supported" ELSE "unsupported"
    [] op = "COMBINE_MEMBERSHIP" ->
         IF stepCount >= 1 THEN "supported" ELSE "unsupported"
    [] op = "ROW_EXPANDED" ->
         IF shape = "records" THEN "supported" ELSE "unsupported"
    [] op = "ROW_EXPLICIT_GROUP" ->
         IF shape = "records" THEN "supported" ELSE "unsupported"
    [] op = "TABLE_SHAPE_PIVOT" ->
         IF ~constructionPresent THEN "supported" ELSE "unsupported"
    [] op = "TABLE_SHAPE_UNPIVOT" ->
         IF ~constructionPresent THEN "supported" ELSE "unsupported"
    [] OTHER -> "unsupported"

(* Mounted-UI availability, including visible-but-disabled controls. *)
UiOffers(op, shape, cols, constructionPresent, stepCount, relatedAnchor) ==
  CASE op = "ADD_FIELD" -> TRUE
    [] op = "ADD_RELATED" -> "related_coding" \in cols
    [] op = "FILTER" -> ScalarCount(cols) >= 1
    [] op = "RELATED_ELIGIBILITY" -> relatedAnchor /\ ScalarCount(cols) >= 1
    [] op = "DERIVE" -> NumericCount(cols) >= 1
    [] op = "GROUP" -> ScalarCount(cols) >= 1
    [] op = "PIVOT" -> ScalarCount(cols) >= 2
    [] op = "CODED_PIVOT" -> "code" \in cols
    [] op = "UNPIVOT" -> ScalarCount(cols) >= 2
    [] op = "EXPAND" ->
         "list_string" \in cols /\ shape = "records" /\ stepCount < 2
    [] op = "RELATED_EXPAND" -> "related_coding" \in cols
    [] op = "COMBINE_APPEND" -> stepCount >= 1
    [] op = "COMBINE_KEY_JOIN" -> stepCount >= 1
    [] op = "COMBINE_MEMBERSHIP" -> stepCount >= 1
    [] op = "ROW_EXPANDED" -> shape = "records"
    [] op = "ROW_EXPLICIT_GROUP" -> shape = "records"
    [] op = "TABLE_SHAPE_PIVOT" -> ~constructionPresent
    [] op = "TABLE_SHAPE_UNPIVOT" -> ~constructionPresent
    [] OTHER -> FALSE

EffectColumns(op, cols) ==
  CASE op = "ADD_FIELD" -> cols \union {"string"}
    [] op = "ADD_RELATED" -> cols \union {"related_coding"}
    [] op = "DERIVE" -> cols \union {"integer"}
    [] op = "GROUP" -> {"string", "integer"}
    [] op = "PIVOT" -> {"string", "integer"}
    [] op = "CODED_PIVOT" -> {"string", "integer"}
    [] op = "UNPIVOT" -> {"string", "integer"}
    [] op = "EXPAND" -> cols \union {"string"}
    [] op = "RELATED_ELIGIBILITY" -> cols
    [] op = "RELATED_EXPAND" -> cols \union {"related_coding"}
    [] op = "COMBINE_APPEND" -> cols \union {"string"}
    [] op = "COMBINE_KEY_JOIN" -> cols \union {"string"}
    [] op = "COMBINE_MEMBERSHIP" -> cols \union {"string"}
    [] op = "ROW_EXPANDED" -> cols
    [] op = "ROW_EXPLICIT_GROUP" -> cols
    [] op = "TABLE_SHAPE_PIVOT" -> {"string", "integer"}
    [] op = "TABLE_SHAPE_UNPIVOT" -> {"string", "integer"}
    [] OTHER -> cols

EffectShape(op) ==
  CASE op = "GROUP" -> "groups"
    [] op = "ROW_EXPLICIT_GROUP" -> "groups"
    [] op = "ROW_EXPANDED" -> "expanded"
    [] op = "EXPAND" -> "expanded"
    [] OTHER -> "records"

VARIABLES
  rowShape, currentColumns, constructionPresent,
  stepPresent, stepOp, stepPrev, stepInputCols, stepCols, stepInputShape,
  restoreCols, restoreShape, candidateSteps, lastStep,
  selectedOp, formValid, relatedAnchor, routedFamily, lastAction

vars == <<
  rowShape, currentColumns, constructionPresent,
  stepPresent, stepOp, stepPrev, stepInputCols, stepCols, stepInputShape,
  restoreCols, restoreShape, candidateSteps, lastStep,
  selectedOp, formValid, relatedAnchor, routedFamily, lastAction
>>

StepCount == Cardinality(candidateSteps)

Init ==
  /\ rowShape = "records"
  /\ currentColumns = BaseColumns
  /\ constructionPresent = FALSE
  /\ stepPresent = [i \in StepIds |-> FALSE]
  /\ stepOp = [i \in StepIds |-> NoOp]
  /\ stepPrev = [i \in StepIds |-> NoStep]
  /\ stepInputCols = [i \in StepIds |-> {}]
  /\ stepCols = [i \in StepIds |-> {}]
  /\ stepInputShape = [i \in StepIds |-> "records"]
  /\ restoreCols = [i \in StepIds |-> BaseColumns]
  /\ restoreShape = [i \in StepIds |-> "records"]
  /\ candidateSteps = {}
  /\ lastStep = NoStep
  /\ selectedOp = NoOp
  /\ formValid = FALSE
  /\ relatedAnchor = TRUE
  /\ routedFamily = "NONE"
  /\ lastAction = "Init"

AuthorStep(op) ==
  /\ op \in AuthorableOps
  /\ StepCount < MaxSteps
  /\ UiOffers(op, rowShape, currentColumns, constructionPresent, StepCount, relatedAnchor)
  /\ StepSupport(op, rowShape, currentColumns, constructionPresent, StepCount, relatedAnchor) = "supported"
  /\ \E id \in StepIds:
       /\ ~stepPresent[id]
       /\ stepPresent' = [stepPresent EXCEPT ![id] = TRUE]
       /\ stepOp' = [stepOp EXCEPT ![id] = op]
       /\ stepPrev' = [stepPrev EXCEPT ![id] = lastStep]
       /\ stepInputCols' = [stepInputCols EXCEPT ![id] = currentColumns]
       /\ stepInputShape' = [stepInputShape EXCEPT ![id] = rowShape]
       /\ stepCols' = [stepCols EXCEPT ![id] = EffectColumns(op, currentColumns)]
       /\ restoreCols' = [restoreCols EXCEPT ![id] = currentColumns]
       /\ restoreShape' = [restoreShape EXCEPT ![id] = rowShape]
       /\ candidateSteps' = candidateSteps \union {id}
       /\ lastStep' = id
       /\ currentColumns' = EffectColumns(op, currentColumns)
       /\ rowShape' = EffectShape(op)
       /\ constructionPresent' = TRUE
       /\ selectedOp' = op
       /\ formValid' = TRUE
       /\ routedFamily' = "NONE"
       /\ relatedAnchor' = relatedAnchor
       /\ lastAction' = "AuthorStep(" \o op \o ")"

UndoLastStep ==
  /\ lastStep # NoStep
  /\ stepPresent' = [stepPresent EXCEPT ![lastStep] = FALSE]
  /\ candidateSteps' = candidateSteps \ {lastStep}
  /\ currentColumns' = restoreCols[lastStep]
  /\ rowShape' = restoreShape[lastStep]
  /\ lastStep' = stepPrev[lastStep]
  /\ constructionPresent' = (candidateSteps' # {})
  /\ lastAction' = "UndoLastStep(" \o stepOp[lastStep] \o ")"
  /\ UNCHANGED <<
       stepOp, stepPrev, stepInputCols, stepCols, stepInputShape,
       restoreCols, restoreShape, selectedOp, formValid, relatedAnchor, routedFamily
     >>

ResetWorkspace ==
  /\ candidateSteps' = {}
  /\ stepPresent' = [i \in StepIds |-> FALSE]
  /\ lastStep' = NoStep
  /\ currentColumns' = BaseColumns
  /\ rowShape' = "records"
  /\ constructionPresent' = FALSE
  /\ selectedOp' = NoOp
  /\ formValid' = FALSE
  /\ routedFamily' = "NONE"
  /\ lastAction' = "ResetWorkspace"
  /\ UNCHANGED <<
       stepOp, stepPrev, stepInputCols, stepCols, stepInputShape,
       restoreCols, restoreShape, relatedAnchor
     >>

RouteSavedStep(id) ==
  /\ id \in StepIds
  /\ stepPresent[id]
  /\ selectedOp' = stepOp[id]
  /\ routedFamily' = EditFamily(stepOp[id])
  /\ lastAction' = "RouteSavedStep(" \o stepOp[id] \o ")"
  /\ UNCHANGED <<
       rowShape, currentColumns, constructionPresent, stepPresent, stepOp, stepPrev,
       stepInputCols, stepCols, stepInputShape, restoreCols, restoreShape,
       candidateSteps, lastStep, formValid, relatedAnchor
     >>

Next ==
  \/ \E op \in AuthorableOps: AuthorStep(op)
  \/ \E id \in StepIds: RouteSavedStep(id)
  \/ UndoLastStep
  \/ ResetWorkspace

Spec == Init /\ [][Next]_vars

Inv_StepsWellFormed ==
  \A id \in StepIds:
    IF stepPresent[id]
    THEN /\ stepInputCols[id] =
              IF stepPrev[id] = NoStep THEN BaseColumns
              ELSE stepCols[stepPrev[id]]
         /\ StepSupport(stepOp[id], stepInputShape[id], stepInputCols[id],
                        FALSE, 1, relatedAnchor) # "unsupported"
    ELSE TRUE

Inv_NoUiOfferedUnsupportedOperation ==
  \A op \in Ops:
    UiOffers(op, rowShape, currentColumns, constructionPresent, StepCount, relatedAnchor) =>
      StepSupport(op, rowShape, currentColumns, constructionPresent,
                  StepCount, relatedAnchor) # "unsupported"

Inv_RoutedEditorMatchesSavedOperation ==
  IF routedFamily = "NONE" THEN TRUE
  ELSE \/ routedFamily = "KEEP_ROWS" /\ selectedOp \in {"FILTER", "RELATED_ELIGIBILITY"}
       \/ routedFamily = "CALCULATE" /\ selectedOp = "DERIVE"
       \/ routedFamily = "RESHAPE" /\ selectedOp \in {"GROUP", "PIVOT", "CODED_PIVOT", "UNPIVOT", "EXPAND"}
       \/ routedFamily = "COMBINE" /\ selectedOp \in {"COMBINE_APPEND", "COMBINE_KEY_JOIN", "COMBINE_MEMBERSHIP"}
       \/ routedFamily = "RELATED" /\ selectedOp \in {"ADD_RELATED", "RELATED_EXPAND"}

TypeOK ==
  /\ rowShape \in Shapes
  /\ currentColumns \in SUBSET ColumnClasses
  /\ constructionPresent \in BOOLEAN
  /\ stepPresent \in [StepIds -> BOOLEAN]
  /\ stepOp \in [StepIds -> OpValues]
  /\ stepPrev \in [StepIds -> LastValues]
  /\ stepInputCols \in [StepIds -> SUBSET ColumnClasses]
  /\ stepCols \in [StepIds -> SUBSET ColumnClasses]
  /\ stepInputShape \in [StepIds -> Shapes]
  /\ restoreCols \in [StepIds -> SUBSET ColumnClasses]
  /\ restoreShape \in [StepIds -> Shapes]
  /\ candidateSteps \in SUBSET StepIds
  /\ lastStep \in LastValues
  /\ selectedOp \in OpValues
  /\ formValid \in BOOLEAN
  /\ relatedAnchor \in BOOLEAN
  /\ routedFamily \in EditFamilies

====
