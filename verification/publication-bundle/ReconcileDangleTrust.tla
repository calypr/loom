------------------------ MODULE ReconcileDangleTrust ------------------------

EXTENDS Naturals, FiniteSets

CONSTANT EXEC,
         ecand,
         AtomicPublish,
         PinAtActivation,
         RecheckBeforeDrop,
         FixedReader

ASSUME Wellformed ==
  /\ EXEC # {}
  /\ ecand \in EXEC
  /\ AtomicPublish \in BOOLEAN
  /\ PinAtActivation \in BOOLEAN
  /\ RecheckBeforeDrop \in BOOLEAN
  /\ FixedReader \in BOOLEAN

CONSTANT RUNNING, PUBLISHED, FAILED

VARIABLES state,
          tableOK,
          pointerWritten,
          candidates,
          checkedAbsent,
          activated

TypeOK ==
  /\ state \in [EXEC -> {RUNNING, PUBLISHED, FAILED}]
  /\ tableOK \in [EXEC -> BOOLEAN]
  /\ pointerWritten \in [EXEC -> BOOLEAN]
  /\ candidates \subseteq EXEC
  /\ checkedAbsent \subseteq candidates
  /\ activated \in BOOLEAN

Init ==
  /\ state = [x \in EXEC |-> IF x = ecand THEN RUNNING ELSE PUBLISHED]
  /\ tableOK = [x \in EXEC |-> TRUE]
  /\ pointerWritten = [x \in EXEC |-> x # ecand]
  /\ candidates = {}
  /\ checkedAbsent = {}
  /\ activated = FALSE

PublishExec ==
  /\ ~AtomicPublish
  /\ state[ecand] = RUNNING
  /\ state' = [state EXCEPT ![ecand] = PUBLISHED]
  /\ UNCHANGED <<tableOK, pointerWritten, candidates, checkedAbsent, activated>>

PublishPointer ==
  /\ ~AtomicPublish
  /\ state[ecand] = PUBLISHED
  /\ ~pointerWritten[ecand]
  /\ pointerWritten' = [pointerWritten EXCEPT ![ecand] = TRUE]
  /\ UNCHANGED <<state, tableOK, candidates, checkedAbsent, activated>>

PublishBoth ==
  /\ AtomicPublish
  /\ state[ecand] = RUNNING
  /\ ~pointerWritten[ecand]
  /\ state' = [state EXCEPT ![ecand] = PUBLISHED]
  /\ pointerWritten' = [pointerWritten EXCEPT ![ecand] = TRUE]
  /\ UNCHANGED <<tableOK, candidates, checkedAbsent, activated>>

Observe ==
  /\ state[ecand] = RUNNING
  /\ ecand \notin candidates
  /\ candidates' = candidates \cup {ecand}
  /\ UNCHANGED <<state, tableOK, pointerWritten, checkedAbsent, activated>>

CheckPointerAbsent ==
  /\ ecand \in candidates
  /\ ~pointerWritten[ecand]
  /\ ecand \notin checkedAbsent
  /\ checkedAbsent' = checkedAbsent \cup {ecand}
  /\ UNCHANGED <<state, tableOK, pointerWritten, candidates, activated>>

Activate ==
  /\ ~activated
  /\ state[ecand] = PUBLISHED
  /\ tableOK[ecand]
  /\ activated' = TRUE
  /\ UNCHANGED <<state, tableOK, pointerWritten, candidates, checkedAbsent>>

DropDemote ==
  /\ ecand \in candidates
  /\ ecand \in checkedAbsent
  /\ ~ (PinAtActivation /\ activated)
  /\ ~ (RecheckBeforeDrop /\ (state[ecand] = PUBLISHED \/ pointerWritten[ecand]))
  /\ state' = [state EXCEPT ![ecand] = FAILED]
  /\ tableOK' = [tableOK EXCEPT ![ecand] = FALSE]
  /\ UNCHANGED <<pointerWritten, candidates, checkedAbsent, activated>>

vars == <<state, tableOK, pointerWritten, candidates, checkedAbsent, activated>>

Next == \/ PublishExec
        \/ PublishPointer
        \/ PublishBoth
        \/ Observe
        \/ CheckPointerAbsent
        \/ Activate
        \/ DropDemote
        \/ UNCHANGED vars

NoDanglingBound ==
  ~activated \/ \A x \in EXEC: state[x] # FAILED /\ tableOK[x]

HealthyDiscovery ==
  ~activated \/ FixedReader \/ \A x \in EXEC: state[x] = PUBLISHED /\ tableOK[x]

Spec == Init /\ [][Next]_vars

=============================================================================
