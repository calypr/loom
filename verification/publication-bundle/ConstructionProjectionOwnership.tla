---------------------- MODULE ConstructionProjectionOwnership ----------------------
(* Bounded bookkeeping model for projection-owner survival (FORMAL-62-U1).
   Quantifies the shipped kept-rule of pruneConstructionSourceProjections
   (internal/explorer/authoringv2/construction_change.go:524-542) against the
   owner-branch acceptance of validatePivotSourceProjection
   (construction_validation.go:339-395): a projection is kept in the shipped
   proposal iff its owner-step survived the change as an owner kind AND some
   surviving step consumes its column. Consumers: release/apply reviewers +
   regression triage (FINDINGS Round 62 §1). No Go test asserts the branch
   reject strings; this model is the reject-direction coverage.

   Source sync authored at 29d2c5b0 (line ranges): owners registry
   construction_change.go:527-530 keeps kinds Pivot|Group|Filter|Derive|Unpivot;
   used-set :531-533; kept rule :536-540; callers :225/:374. Validation switch
   construction_validation.go:348-377 (pivot/group consumes inputColumnIDs
   :349-352; filter nil-guard + Filter.ColumnID equality :353-356; derive
   nil-guard + COLUMN operand :357-360; unpivot nil-guard + melt membership
   :361-371; default reject :372-374); owner-exists :343-346. Consumption sets
   Operation.inputColumnIDs construction_change.go:839-885.

   Bounds (honest scope): Owners = {pivot,filter,derive,unpivot,sort};
   "group" shares the pivot/group case label (unowned-legacy group path
   validateLegacyGroupSourceProjection out of bound); one projection per
   owner with distinct columns; owner at step index 0 so the carry-prefix
   loop :378-393 is unmodeled; nil payloads subsume into a false consumed
   bit (explicit nil-guards :354/:358/:363 are the reject string, same
   accept-decision). *)

EXTENDS Naturals, TLC

CONSTANTS FPruneDropsSurviving,    \* TRUE: prune drops a projection whose
                                   \*   owner survived and is used (the
                                   \*   FORMAL-62-U1 named wrong-fix shape)
          FPruneKeepsOrphan,       \* TRUE: kept rule ignores the owners[]
                                   \*     registry (projection of a removed
                                   \*     owner survives)
          FPruneIgnoresUsed,       \* TRUE: kept rule ignores the used[] set
          FBranchIgnoresPivotGroup, \* TRUE: case pivot/group vacuous (:349-352)
          FBranchIgnoresFilter,    \* TRUE: case filter vacuous (:353-356)
          FBranchIgnoresDerive,    \* TRUE: case derive vacuous (:357-360)
          FBranchIgnoresUnpivot,   \* TRUE: case unpivot vacuous (:361-371)
          FBranchIgnoresDefault    \* TRUE: default branch vacuous (:372-374)

ASSUME /\ FPruneDropsSurviving \in BOOLEAN
       /\ FPruneKeepsOrphan \in BOOLEAN
       /\ FPruneIgnoresUsed \in BOOLEAN
       /\ FBranchIgnoresPivotGroup \in BOOLEAN
       /\ FBranchIgnoresFilter \in BOOLEAN
       /\ FBranchIgnoresDerive \in BOOLEAN
       /\ FBranchIgnoresUnpivot \in BOOLEAN
       /\ FBranchIgnoresDefault \in BOOLEAN

Owners       == {"pivot", "filter", "derive", "unpivot", "sort"}
OwnerKinds   == {"pivot", "filter", "derive", "unpivot"}   \* construction_change.go:528-530

VARIABLES stepPresent,   \* [Owners -> BOOLEAN]: owner step survived the change
          projPresent,   \* [Owners -> BOOLEAN]: candidate carries the projection
          consumed,      \* [Owners -> BOOLEAN]: owner consumes column (kind rule)
          crossUsed,     \* [Owners -> BOOLEAN]: another surviving step consumes it
          kept,          \* [Owners -> BOOLEAN]: prune kept the projection
          result,        \* "choosing" | "none" | "persisted" | "rejected"
          chosen         \* owners whose inputs are fixed

vars == <<stepPresent, projPresent, consumed, crossUsed, kept, result, chosen>>

\* Global used[] set: owner consumes its column, or any other surviving step
\* does (construction_change.go:531-533 abstracted by crossUse bits).
usedBy(o) == (stepPresent[o] /\ consumed[o]) \/ crossUsed[o]

\* Owners[] registry lookup: a step that survived with an owner-kind is in
\* owners; removed steps and the non-owner kind are not (:527-530 / :537).
ownerMap(o) == stepPresent[o] /\ o \in OwnerKinds

keepRule(o) ==
  IF projPresent[o]
  THEN /\ usedBy(o) \/ FPruneIgnoresUsed
       /\ ownerMap(o) \/ FPruneKeepsOrphan
       /\ ~ (FPruneDropsSurviving /\ usedBy(o) /\ ownerMap(o))
  ELSE FALSE

\* Branch acceptance per case label; kept-but-unaccepted rejects the proposal.
branchAccepts(o) ==
  IF ~ stepPresent[o] THEN FALSE                 \* owner step does not exist :343-346
  ELSE IF o = "pivot" \/ o = "group"
    THEN consumed[o] \/ FBranchIgnoresPivotGroup  \* :349-352
  ELSE IF o = "filter"
    THEN consumed[o] \/ FBranchIgnoresFilter      \* :353-356
  ELSE IF o = "derive"
    THEN consumed[o] \/ FBranchIgnoresDerive      \* :357-360
  ELSE IF o = "unpivot"
    THEN consumed[o] \/ FBranchIgnoresUnpivot     \* :361-371
  ELSE FBranchIgnoresDefault                      \* :372-374

Init ==
  /\ stepPresent = [o \in Owners |-> FALSE]
  /\ projPresent = [o \in Owners |-> FALSE]
  /\ consumed    = [o \in Owners |-> FALSE]
  /\ crossUsed   = [o \in Owners |-> FALSE]
  /\ kept        = [o \in Owners |-> FALSE]
  /\ result = "choosing"
  /\ chosen = {}

\* Choices are quantified at the action level (house idiom, mirroring
\* ConstructionPrepareLanes Propose): \E on an EXCEPT right-hand side does
\* not branch under this tla2tools build (observed: 34-state collapse).
BitChoices == [ {"step", "proj", "cons", "cross"} -> BOOLEAN ]

ChooseOwner(o, c) ==
  /\ o \in Owners \ chosen
  /\ stepPresent' = [stepPresent EXCEPT ![o] = c.step]
  /\ projPresent' = [projPresent EXCEPT ![o] = c.proj]
  /\ consumed'    = [consumed EXCEPT ![o] = c.cons]
  /\ crossUsed'   = [crossUsed EXCEPT ![o] = c.cross]
  /\ UNCHANGED <<kept, result>>
  /\ chosen' = chosen \cup {o}

Prune ==
  /\ chosen = Owners
  /\ result = "choosing"
  /\ kept' = [o \in Owners |-> keepRule(o)]
  /\ result' = "none"
  /\ UNCHANGED <<stepPresent, projPresent, consumed, crossUsed, chosen>>

Validate ==
  /\ result = "none"
  /\ result' = IF \E o \in Owners: kept[o] /\ ~ branchAccepts(o)
               THEN "rejected"
               ELSE "persisted"
  /\ UNCHANGED <<stepPresent, projPresent, consumed, crossUsed, kept, chosen>>

Next == (\E o \in Owners, c \in BitChoices: ChooseOwner(o, c)) \/ Prune \/ Validate

Spec == Init /\ [][Next]_vars

TypeOK ==
  /\ stepPresent \in [Owners -> BOOLEAN]
  /\ projPresent \in [Owners -> BOOLEAN]
  /\ consumed \in [Owners -> BOOLEAN]
  /\ crossUsed \in [Owners -> BOOLEAN]
  /\ kept \in [Owners -> BOOLEAN]
  /\ result \in {"choosing", "none", "persisted", "rejected"}
  /\ chosen \subseteq Owners

\* THE named promise: kept in the shipped proposal iff the owner survived
\* prune as an owner kind AND the column is used. One direction failing =
\* orphan shipped (FPruneKeepsOrphan, FPruneIgnoresUsed); the other =
\* legitimately-owned projection silently dropped (FPruneDropsSurviving).
KeepIffOwnerSurvived ==
  result = "choosing"
    \/ \A o \in Owners:
         kept[o] <=> (projPresent[o] /\ usedBy(o) /\ ownerMap(o))

\* Shipped projections are consumed by existing owners: nothing reaches
\* Validate-persisted that its owner-kind branch would reject.
ShippedRequiresConsumption ==
  result # "persisted"
    \/ \A o \in Owners: kept[o] => (stepPresent[o] /\ consumed[o])

=============================================================================
