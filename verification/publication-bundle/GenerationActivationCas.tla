----------------------- MODULE GenerationActivationCas -----------------------

EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS Services, Generations, CasEnforced

ASSUME
  /\ Services \subseteq STRING
  /\ Cardinality(Services) = 2
  /\ Generations \subseteq Nat
  /\ Cardinality(Generations) >= 2
  /\ CasEnforced \in BOOLEAN

NOT_YET == 99
GenA == 1
GenB == 2

VARIABLES
  ptr,
  view,
  decided,
  activated,
  errored,
  blinded

firstService == CHOOSE s \in Services : TRUE
target(s) == IF s = firstService THEN GenA ELSE GenB

TypeInit ==
  /\ ptr = 0
  /\ view \in [Services -> {0}]
  /\ decided \in [Services -> BOOLEAN]
  /\ activated \in [Services -> {NOT_YET}]
  /\ errored \in [Services -> BOOLEAN]
  /\ blinded = [s \in Services |-> FALSE]

Read(s) ==
  /\ activated[s] = NOT_YET
  /\ decided[s] = FALSE
  /\ decided' = [decided EXCEPT ![s] = TRUE]
  /\ view' = [view EXCEPT ![s] = ptr]
  /\ UNCHANGED <<ptr, activated, errored, blinded>>

Activate(s) ==
  /\ activated[s] = NOT_YET
  /\ decided[s] = TRUE
  /\ IF CasEnforced
       THEN IF view[s] = ptr
              THEN /\ activated' = [activated EXCEPT ![s] = target(s)]
                   /\ ptr' = target(s)
                   /\ UNCHANGED <<view, decided, errored, blinded>>
              ELSE /\ errored' = [errored EXCEPT ![s] = TRUE]
                   /\ UNCHANGED <<ptr, activated, view, decided, blinded>>
       ELSE /\ activated' = [activated EXCEPT ![s] = target(s)]
            /\ ptr' = target(s)
            /\ blinded' = [blinded EXCEPT ![s] = blinded[s] \/ (view[s] # ptr)]
            /\ UNCHANGED <<view, decided, errored>>

Retry(s) ==
  /\ errored[s]
  /\ decided' = [decided EXCEPT ![s] = FALSE]
  /\ UNCHANGED <<ptr, view, activated, errored, blinded>>

Loop == UNCHANGED <<ptr, view, decided, activated, errored, blinded>>

Next ==
  \/ \E s \in Services : Read(s)
  \/ \E s \in Services : Activate(s)
  \/ \E s \in Services : Retry(s)
  \/ Loop

Init == TypeInit

stvars == <<ptr, view, decided, activated, errored, blinded>>

NoBlindCommit ==
  \A s \in Services : \lnot blinded[s]

FairSpec ==
  Init /\ [][Next]_stvars
  /\ WF_stvars(\E s \in Services : Read(s))
  /\ WF_stvars(\E s \in Services : Activate(s))
  /\ WF_stvars(\E s \in Services : Retry(s))
=============================================================================
