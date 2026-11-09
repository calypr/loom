--------------------------- MODULE PointerOrphanLifecycle ---------------------------
(* Formal witness for candidate F-B19: PUBLISHED executions orphaned by a newer
   publish under the same pointer name were never garbage-collected, because the
   pre-repair reconcile sweep covered only {RUNNING, VALIDATING, FAILED}
   (pre-FIX clickhouse/bundle_store.go) and removal otherwise requires the owner
   lease (arango/bundle_registry.go REMOVE lease-only). Controls: a recurring
   janitor converges, and only a janitor that respects active-release bindings
   is safe (else it re-creates the F-B12-shaped bound-data loss).

   SOURCE SYNC (Round 32, Repair-4 SHIPPED): clickhouse/bundle_store.go
   Reconcile now appends BundlePublished to the sweep states ONLY when an
   ActiveReleaseReader is wired (server.go production wiring); pre-claim
   filter skips pointer-named / active-release-bound PUBLISHED rows WITHOUT
   claiming; destruction still requires the post-claim pointer + binding
   re-checks (RecheckBeforeReclaim = TRUE arm) with fail-closed reads, and a
   cleanup-lease+pin claim. Shipped behavior = this model's m-po-fixed
   constant set (JanitorPresent, JanitorChecksBindings, RecheckBeforeReclaim
   all TRUE). m-po-bug (no janitor) remains the archived F-B19 witness and
   the pre-fix e2e behavior; m-po-janitor-blind is why PUBLISHED is NOT
   enumerated without a bound reader; m-po-reclaim-blind underpins the
   Repair-1b drop-path veto. *)

EXTENDS Naturals, FiniteSets

CONSTANTS MaxExecs, JanitorPresent, JanitorChecksBindings, RecheckBeforeReclaim

ASSUME MaxExecs \in Nat /\ MaxExecs >= 2

VARIABLES
  nextKey,        \* monotone key supply; each publish mints a fresh Key
  state,          \* [Key -> {"RUNNING","VALIDATING","FAILED","PUBLISHED","GONE"}]
  pointer,        \* Key named by the (single) pointer row (always in Keys)
  boundKeys,      \* set of Keys referenced by active release bindings
  leases          \* set of Keys whose owner lease is still live (REMOVE requires it)

Keys == 1..MaxExecs
LiveStates == {"RUNNING","VALIDATING","FAILED","PUBLISHED"}

stvars == <<nextKey, state, pointer, boundKeys, leases>>

TypeInit ==
  /\ nextKey = 1
  /\ state \in [Keys -> LiveStates \cup {"GONE"}]
  \* model starts with one already-superseded PUBLISHED orphan (F-B19 regime):
  \* key 1 PUBLISHED, pointer names key 2, nothing bound.
  /\ state[1] = "PUBLISHED"
  /\ state[2] = "PUBLISHED"
  /\ pointer = 2
  /\ boundKeys = {}
  /\ leases = {}   \* prior owners' leases all expired; orphans are lease-less

(* PublishExecution leg: fresh Key, both writes land (single-commit arm).
   The old PUBLISHED exec keeps existing; the pointer row is overwritten. *)
Publish(e) ==
  /\ nextKey = e
  /\ e <= MaxExecs
  /\ state' = [state EXCEPT ![e] = "PUBLISHED"]
  /\ pointer' = e
  /\ nextKey' = e + 1
  /\ UNCHANGED boundKeys
  /\ leases' = leases \cup {e}   \* publisher holds a live lease on its fresh exec

(* Active release re-point: binds whatever the pointer names now; the
   previously bound exec may become orphaned by this action. *)
Rebind ==
  /\ state[pointer] = "PUBLISHED"
  /\ boundKeys' = {pointer}
  /\ UNCHANGED <<nextKey, state, pointer, leases>>

(* Shipped reconcile sweep: touches only the three pre-publish states. *)
ShippedSweep(e) ==
  /\ state[e] \in {"RUNNING","VALIDATING","FAILED"}
  /\ state' = [state EXCEPT ![e] = "GONE"]
  /\ UNCHANGED <<nextKey, pointer, boundKeys, leases>>

(* Owner-lease removal: THE ONLY path to GONE for a PUBLISHED exec today,
   gated on the live lease (arango/bundle_registry.go REMOVE lease-only). *)
LeaseRemove(e) ==
  /\ state[e] # "GONE"
  /\ e \in leases
  /\ pointer # e        \* shipped pointer guard (bundle_store.go:1068-1075 analog)
  /\ IF RecheckBeforeReclaim THEN e \notin boundKeys ELSE TRUE   \* fix menu item 1
  /\ state' = [state EXCEPT ![e] = "GONE"]
  /\ leases' = leases \ {e}
  /\ UNCHANGED <<nextKey, pointer, boundKeys>>

LeaseExpire(e) ==
  /\ e \in leases
  /\ leases' = leases \ {e}
  /\ UNCHANGED <<nextKey, state, pointer, boundKeys>>

(* Proposed control: recurring janitor for PUBLISHED orphans. *)
Orphan(e) ==
  /\ state[e] = "PUBLISHED"
  /\ pointer # e
  /\ e \notin boundKeys

Janitor(e) ==
  /\ JanitorPresent
  /\ Orphan(e)
  /\ IF JanitorChecksBindings THEN TRUE ELSE TRUE  \* binding check modeled below
  /\ state' = [state EXCEPT ![e] = "GONE"]
  /\ UNCHANGED <<nextKey, pointer, boundKeys, leases>>

(* Unsafe janitor arm: ignores bindings, may reap a bound exec. *)
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

(* F-B19: every orphan is eventually collected. Shipped sweep never touches
   PUBLISHED, so this must be violated when no janitor exists. *)
(* Only PERMANENTLY orphaned execs are the harm: transient orphanhood that a
   later rebind rescues is legitimate product behavior (release re-pointing). *)
OrphanGC ==
  \A e \in Keys : []( [](Orphan(e)) => <>(state[e] = "GONE") )

(* Safety demanded of the fix: never reap what an active release binds. *)
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
