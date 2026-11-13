----------------------- MODULE GenerationActivationCas -----------------------
(* Adversarial witness for confirmed bug F-B17: the shared active_generation
   pointer is flipped by manifest_lifecycle.go activateAQL with NO value/
   revision/lease compare (UPDATE active WITH {dataset, manifestKey} OPTIONS
   {ignoreRevs:false, mergeObjects:false} at :334-335). The read-then-act
   pre-check ResolveActiveManifest at :198-204 is a separate round trip whose
   result the write never re-reads. The bulk-load caller
   (internal/api/bulk/load/service.go :206 resolve -> :210 IsStaged -> :213
   Activate) adds no protection. User harm: Activate documents/promises that a
   nil return leaves the caller's generation active; when two services run the
   pattern concurrently for different generations, the loser's activation is
   silently discarded yet it still observes err=nil.

   This model establishes the promise violation is a MODEL-level fact of the
   shipped shape and that the quoted fix shape (compare-against-observed-value
   before patch, cf. release_store.go:166-167) closes it. The e2e
   (parent run 98155374) is the real-Arango confirmation; this is the formal
   control arm, NOT a substitute for it. *)

EXTENDS Naturals, FiniteSets, Sequences

CONSTANTS Services, Generations, CasEnforced

ASSUME
  /\ Services \subseteq STRING
  /\ Cardinality(Services) = 2
  /\ Generations \subseteq Nat
  /\ Cardinality(Generations) >= 2
  /\ CasEnforced \in BOOLEAN

(* Two distinct target generations, one per service; NOT_YET is the
   un-activated sentinel (distinct from every target and from 0). *)
NOT_YET == 99
GenA == 1
GenB == 2

VARIABLES
  ptr,        \* the generation the shared active_generation pointer names (0 == none)
  view,       \* [service -> generation observed at the :206 resolve step]
  decided,    \* [service -> TRUE once it has resolved (single decision per wave)]
  activated,  \* [service -> generation its Activate committed, or NOT_YET]
  errored,    \* [service -> TRUE if its Activate surfaced a conflict instead]
  blinded     \* ghost: [service -> TRUE once it COMMITTED over a pointer value
                       \*       it never observed — the F-B17 harm predicate]

(* Deterministic distinct target per service: the first CHOOSE-named service
   gets GenA, the other gets GenB. The identity of "first" is irrelevant to the
   property (only distinctness + both-successes symmetry matter). *)
firstService == CHOOSE s \in Services : TRUE
target(s) == IF s = firstService THEN GenA ELSE GenB

TypeInit ==
  /\ ptr = 0
  /\ view \in [Services -> {0}]
  /\ decided \in [Services -> BOOLEAN]
  /\ activated \in [Services -> {NOT_YET}]
  /\ errored \in [Services -> BOOLEAN]
  /\ blinded = [s \in Services |-> FALSE]

(* :206 resolve step. Latches a single decision per service; the value the
   later write is (in the fix shape) supposed to still match. *)
Read(s) ==
  /\ activated[s] = NOT_YET
  /\ decided[s] = FALSE
  /\ decided' = [decided EXCEPT ![s] = TRUE]
  /\ view' = [view EXCEPT ![s] = ptr]
  /\ UNCHANGED <<ptr, activated, errored, blinded>>

(* :213 Activate. Fix shape (CasEnforced) re-checks that the pointer still
   equals what it observed at :206; the shipped shape (blanket else) overwrites
   unconditionally. *)
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
            \* ghost faithful to the shipped AQL: the prior pointer value is
            \* compared against NOTHING; blinded records whether the committer
            \* had ever observed it.
            /\ blinded' = [blinded EXCEPT ![s] = blinded[s] \/ (view[s] # ptr)]
            /\ UNCHANGED <<view, decided, errored>>

(* A conflicted caller may re-gate (fresh single observation) — modeling the
   honest retry loop; a fresh gate is a lawful last-writer-wins read. *)
Retry(s) ==
  /\ errored[s]
  /\ decided' = [decided EXCEPT ![s] = FALSE]
  /\ UNCHANGED <<ptr, view, activated, errored, blinded>>

(* explicit loopback so no state is terminal (stuttering made first-class) *)
Loop == UNCHANGED <<ptr, view, decided, activated, errored, blinded>>

Next ==
  \/ \E s \in Services : Read(s)
  \/ \E s \in Services : Activate(s)
  \/ \E s \in Services : Retry(s)
  \/ Loop

Init == TypeInit

stvars == <<ptr, view, decided, activated, errored, blinded>>

(* F-B17 harm predicate: no service may COMMIT an activation over a pointer
   value it never observed at its gate. Shipped blind activateAQL violates
   (R1 reads 0, R2 reads 0, R1 commits => ptr moves; R2's commit over the
   moved pointer was never gated on it). The expected-value CAS shape can only
   commit when view[s] = ptr, so blinded is never set — including legitimate
   late-gate last-writer-wins, which stays lawful. *)
NoBlindCommit ==
  \A s \in Services : \lnot blinded[s]

FairSpec ==
  Init /\ [][Next]_stvars
  /\ WF_stvars(\E s \in Services : Read(s))
  /\ WF_stvars(\E s \in Services : Activate(s))
  /\ WF_stvars(\E s \in Services : Retry(s))
=============================================================================
