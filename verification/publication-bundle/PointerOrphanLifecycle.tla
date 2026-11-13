--------------------------- MODULE PointerOrphanLifecycle ---------------------------

EXTENDS Naturals, FiniteSets

CONSTANTS MaxExecs, JanitorPresent, JanitorChecksBindings, RecheckBeforeReclaim

ASSUME MaxExecs \in Nat /\ MaxExecs >= 2

VARIABLES
  nextKey,
  state,
  pointer,
  boundKeys,
  leases

Keys == 1..MaxExecs
LiveStates == {"RUNNING","VALIDATING","FAILED","PUBLISHED"}

stvars == <<nextKey, state, pointer, boundKeys, leases>>

TypeInit ==
  /\ nextKey = 1
  /\ state \in [Keys -> LiveStates \cup {"GONE"}]
  /\ state[1] = "PUBLISHED"
  /\ state[2] = "PUBLISHED"
  /\ pointer = 2
  /\ boundKeys = {}
  /\ leases = {}

Publish(e) ==
  /\ nextKey = e
  /\ e <= MaxExecs
  /\ state' = [state EXCEPT ![e] = "PUBLISHED"]
  /\ pointer' = e
  /\ nextKey' = e + 1
  /\ UNCHANGED boundKeys
  /\ leases' = leases \cup {e}

Rebind ==
  /\ state[pointer] = "PUBLISHED"
  /\ boundKeys' = {pointer}
  /\ UNCHANGED <<nextKey, state, pointer, leases>>

ShippedSweep(e) ==
  /\ state[e] \in {"RUNNING","VALIDATING","FAILED"}
  /\ state' = [state EXCEPT ![e] = "GONE"]
  /\ UNCHANGED <<nextKey, pointer, boundKeys, leases>>

LeaseRemove(e) ==
  /\ state[e] # "GONE"
  /\ e \in leases
  /\ pointer # e
  /\ IF RecheckBeforeReclaim THEN e \notin boundKeys ELSE TRUE
  /\ state' = [state EXCEPT ![e] = "GONE"]
  /\ leases' = leases \ {e}
  /\ UNCHANGED <<nextKey, pointer, boundKeys>>

LeaseExpire(e) ==
  /\ e \in leases
  /\ leases' = leases \ {e}
  /\ UNCHANGED <<nextKey, state, pointer, boundKeys>>

Orphan(e) ==
  /\ state[e] = "PUBLISHED"
  /\ pointer # e
  /\ e \notin boundKeys

Janitor(e) ==
  /\ JanitorPresent
  /\ Orphan(e)
  /\ IF JanitorChecksBindings THEN TRUE ELSE TRUE
  /\ state' = [state EXCEPT ![e] = "GONE"]
  /\ UNCHANGED <<nextKey, pointer, boundKeys, leases>>

JanitorBlind(e) ==
  /\ JanitorPresent
  /\ \lnot JanitorChecksBindings
  /\ IF RecheckBeforeReclaim THEN e \notin boundKeys ELSE TRUE
  /\ state[e] = "PUBLISHED"
  /\ pointer # e
  /\ state' = [state EXCEPT ![e] = "GONE"]
  /\ UNCHANGED <<nextKey, pointer, boundKeys, leases>>

Next ==
  \/ \E e \in Keys : Publish(e)
  \/ Rebind
  \/ \E e \in Keys : ShippedSweep(e)
  \/ \E e \in Keys : LeaseRemove(e)
  \/ \E e \in Keys : LeaseExpire(e)
  \/ \E e \in Keys : Janitor(e)
  \/ \E e \in Keys : JanitorBlind(e)

Init == TypeInit

OrphanGC ==
  \A e \in Keys : []( [](Orphan(e)) => <>(state[e] = "GONE") )

NoBoundDeleted ==
  \A e \in boundKeys : state[e] # "GONE"

FairSpec ==
  Init /\ [][Next]_stvars
  /\ WF_stvars(\E e \in Keys : Publish(e))
  /\ WF_stvars(\E e \in Keys : ShippedSweep(e))
  /\ WF_stvars(\E e \in Keys : LeaseRemove(e))
  /\ WF_stvars(\E e \in Keys : LeaseExpire(e))
  /\ WF_stvars(\E e \in Keys : Janitor(e))
  /\ WF_stvars(\E e \in Keys : JanitorBlind(e))
=============================================================================
