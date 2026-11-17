---------------------- MODULE ConstructionProjectionOwnership ----------------------

EXTENDS Naturals, TLC

CONSTANTS FPruneDropsSurviving,
          FPruneKeepsOrphan,
          FPruneIgnoresUsed,
          FBranchIgnoresPivotGroup,
          FBranchIgnoresFilter,
          FBranchIgnoresDerive,
          FBranchIgnoresUnpivot,
          FBranchIgnoresDefault

ASSUME /\ FPruneDropsSurviving \in BOOLEAN
       /\ FPruneKeepsOrphan \in BOOLEAN
       /\ FPruneIgnoresUsed \in BOOLEAN
       /\ FBranchIgnoresPivotGroup \in BOOLEAN
       /\ FBranchIgnoresFilter \in BOOLEAN
       /\ FBranchIgnoresDerive \in BOOLEAN
       /\ FBranchIgnoresUnpivot \in BOOLEAN
       /\ FBranchIgnoresDefault \in BOOLEAN

Owners       == {"pivot", "filter", "derive", "unpivot", "sort"}
OwnerKinds   == {"pivot", "filter", "derive", "unpivot"}

VARIABLES stepPresent,
          projPresent,
          consumed,
          crossUsed,
          kept,
          result,
          chosen

vars == <<stepPresent, projPresent, consumed, crossUsed, kept, result, chosen>>

usedBy(o) == (stepPresent[o] /\ consumed[o]) \/ crossUsed[o]

ownerMap(o) == stepPresent[o] /\ o \in OwnerKinds

keepRule(o) ==
  IF projPresent[o]
  THEN /\ usedBy(o) \/ FPruneIgnoresUsed
       /\ ownerMap(o) \/ FPruneKeepsOrphan
       /\ ~ (FPruneDropsSurviving /\ usedBy(o) /\ ownerMap(o))
  ELSE FALSE

branchAccepts(o) ==
  IF ~ stepPresent[o] THEN FALSE
  ELSE IF o = "pivot" \/ o = "group"
    THEN consumed[o] \/ FBranchIgnoresPivotGroup
  ELSE IF o = "filter"
    THEN consumed[o] \/ FBranchIgnoresFilter
  ELSE IF o = "derive"
    THEN consumed[o] \/ FBranchIgnoresDerive
  ELSE IF o = "unpivot"
    THEN consumed[o] \/ FBranchIgnoresUnpivot
  ELSE FBranchIgnoresDefault

Init ==
  /\ stepPresent = [o \in Owners |-> FALSE]
  /\ projPresent = [o \in Owners |-> FALSE]
  /\ consumed    = [o \in Owners |-> FALSE]
  /\ crossUsed   = [o \in Owners |-> FALSE]
  /\ kept        = [o \in Owners |-> FALSE]
  /\ result = "choosing"
  /\ chosen = {}

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

KeepIffOwnerSurvived ==
  result = "choosing"
    \/ \A o \in Owners:
         kept[o] <=> (projPresent[o] /\ usedBy(o) /\ ownerMap(o))

ShippedRequiresConsumption ==
  result # "persisted"
    \/ \A o \in Owners: kept[o] => (stepPresent[o] /\ consumed[o])

=============================================================================
