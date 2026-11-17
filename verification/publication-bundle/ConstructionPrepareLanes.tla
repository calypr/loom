------------------------- MODULE ConstructionPrepareLanes -------------------------

EXTENDS Naturals, TLC

CONSTANTS FHelperGateIgnoresCleared,
          FSetBranchGateIgnoresCleared,
          FSetBranchPersistsUnconsumed,
          FSharedClearedGate

ASSUME /\ FHelperGateIgnoresCleared \in BOOLEAN
       /\ FSetBranchGateIgnoresCleared \in BOOLEAN
       /\ FSetBranchPersistsUnconsumed \in BOOLEAN
       /\ FSharedClearedGate \in BOOLEAN

Lanes  == {"pivot", "filter", "derive", "unpivot"}
Carries == {"Untouched", "Omitted", "Cleared", "Replaced"}
States == {"None", "Intact"}

VARIABLES durable,
          prev,
          picks,
          lastCarried,
          result

vars == <<durable, prev, picks, lastCarried, result>>

clearedPred(base, carry, o) == /\ base[o] = "Intact"
                               /\ carry[o] = "Cleared"
anyCleared(base, carry) == \E e \in Lanes: clearedPred(base, carry, e)

helperDomain(L, o)   == o = "pivot" /\ L # "pivot"
setBranchSite(L)     == L \in {"pivot", "filter"}
frozenSite(L)        == L \in {"derive", "unpivot"}

faultRestores(L, base, carry, o) ==
  IF o # L /\ clearedPred(base, carry, o)
  THEN \/ /\ helperDomain(L, o) /\ FHelperGateIgnoresCleared
       \/ /\ setBranchSite(L) /\ FSetBranchGateIgnoresCleared
       \/ /\ frozenSite(L) /\ FSetBranchPersistsUnconsumed
       \/ FALSE
  ELSE FALSE

proposalError(L, base, carry) ==
  \E o \in Lanes: faultRestores(L, base, carry, o)
                 /\ ~(frozenSite(L) /\ FSetBranchPersistsUnconsumed)

newState(L, base, carry, o) ==
  IF o = L
    THEN IF carry[o] = "Replaced" THEN "Intact"
         ELSE IF carry[o] = "Cleared" THEN "None"
         ELSE base[o]
    ELSE IF base[o] = "Intact"
      THEN IF carry[o] = "Cleared"
        THEN IF faultRestores(L, base, carry, o)
             THEN IF frozenSite(L) /\ FSetBranchPersistsUnconsumed
                  THEN "Intact"
                  ELSE base[o]
             ELSE "None"
        ELSE IF carry[o] = "Omitted"
          THEN IF FSharedClearedGate /\ anyCleared(base, carry)
               THEN "None"
               ELSE "Intact"
          ELSE IF carry[o] = "Replaced" THEN "Intact"
          ELSE base[o]
      ELSE IF carry[o] = "Replaced" THEN "Intact"
      ELSE "None"

Init ==
  /\ \E d \in [Lanes -> States]: /\ durable = d
                                 /\ prev = d
  /\ picks = {}
  /\ lastCarried = [o \in Lanes |-> "never"]
  /\ result = "started"

Propose(L, carry) ==
  /\ L \in Lanes \ picks
  /\ carry \in [Lanes -> Carries]
  /\ picks' = picks \cup {L}
  /\ prev' = durable
  /\ lastCarried' = carry
  /\ IF proposalError(L, durable, carry)
       THEN /\ durable' = durable
            /\ result' = "error"
       ELSE /\ durable' = [o \in Lanes |-> newState(L, durable, carry, o)]
            /\ result' = "persisted"

Next == \E L \in Lanes, carry \in [Lanes -> Carries]: Propose(L, carry)

Spec == Init /\ [][Next]_vars

TypeOK ==
  /\ durable \in [Lanes -> States]
  /\ prev \in [Lanes -> States]
  /\ picks \subseteq Lanes
  /\ lastCarried \in [Lanes -> (Carries \cup {"never"})]
  /\ result \in {"started", "persisted", "error"}

ClearedStaysCleared ==
  \A o \in Lanes: lastCarried[o] = "Cleared" => /\ result = "persisted"
                                                 /\ durable[o] = "None"

NoFalseRestore ==
  \A o \in Lanes: /\ durable[o] = "Intact"
                  /\ prev[o] = "None"
               => lastCarried[o] = "Replaced"

UntouchedOwnerSurvival ==
  \A o \in Lanes: /\ prev[o] = "Intact"
                  /\ lastCarried[o] \in {"Untouched", "Omitted"}
                  /\ result = "persisted"
               => durable[o] = "Intact"

OwnerIsolation ==
  \A o \in Lanes: durable[o] # prev[o]
               => lastCarried[o] \in {"Cleared", "Replaced"}

=============================================================================
