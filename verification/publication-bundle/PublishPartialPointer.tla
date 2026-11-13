--------------------------- MODULE PublishPartialPointer ---------------------------
EXTENDS Integers, TLC

\* Formal control for the F-B16 mechanism class. Pre-fix (dc9016bc),
\* PublishExecution committed the execution doc and the pointer doc in ONE
\* non-transactional multi-statement AQL (bundle_registry.go:292-296 @ dc9016bc);
\* a mid-query abort could leave the execution PUBLISHED while the pointer
\* never names it. SOURCE SYNC (round 26 repair): both legs now run inside one
\* Arango transaction (WithTransaction over executions+pointers+leases), so
\* AtomicPublish = TRUE is the shipped shape and m-ppp-atomic is the
\* current-code baseline; AtomicPublish = FALSE (m-ppp-bug) is retained as the
\* pre-fix fault control and models legacy rows created before the repair.
\* Production fallbacks then close
\* the tx uncertain (bundle_store.go:818-821), idempotent replays return the
\* published result without repair (beginBundle :137-140; Commit :762-764), and
\* reconcile only sweeps RUNNING/VALIDATING/FAILED (:1043). Two product
\* "current" surfaces permanently disagree: the pointer-equality bulk-load gate
\* (bulk/load/service.go:199-204) vs the pointer-blind name-latest selector
\* (bundle_registry.go:174-182).

\* CONTROLS:
\*   AtomicPublish        true => commit publishes state and pointer together.
\*   RepairOnIdempotent   true => an idempotent replay of the same identity
\*                          re-points the pointer for a PUBLISHED execution.
\*   SweepPublished       true => reconcile enumerates PUBLISHED executions and
\*                          repairs pointer-inconsistent ones (counterfactual
\*                          fix; today's sweep excludes PUBLISHED, :1043).

CONSTANT EXEC
CONSTANT AtomicPublish
CONSTANT RepairOnIdempotent
CONSTANT SweepPublished

VARIABLES
  state,        \* EXEC -> { "RUNNING", "PUBLISHED" }
  pointer,      \* EXEC -> { NIL, PRESENT }
  swept         \* EXEC -> BOOLEAN

vars == <<state, pointer, swept>>

Nil == "NIL"
Present == "PRESENT"

TypeOK ==
  /\ state \in [EXEC -> {"RUNNING", "PUBLISHED"}]
  /\ pointer \in [EXEC -> {Nil, Present}]
  /\ swept \in [EXEC -> BOOLEAN]

Init ==
  /\ state = [e \in EXEC |-> "RUNNING"]
  /\ pointer = [e \in EXEC |-> Nil]
  /\ swept = [e \in EXEC |-> FALSE]

\* The PublishExecution AQL as ONE action with two non-deterministic outcomes.
\* Normal leg lands both docs. Partial leg (only when the query is NOT atomic)
\* applies the exec UPDATE (292) then aborts before the pointer UPSERT lands
\* (293-296). The pointer NEVER lands independently of the commit: a retry goes
\* through the idempotent path, not the AQL.
Commit(e) ==
  /\ state[e] = "RUNNING"
  /\ pointer[e] = Nil
  /\ \/ /\ state' = [state EXCEPT ![e] = "PUBLISHED"]
       /\ pointer' = [pointer EXCEPT ![e] = Present]
     \/ /\ AtomicPublish = FALSE
       /\ state' = [state EXCEPT ![e] = "PUBLISHED"]
       /\ UNCHANGED pointer
  /\ UNCHANGED swept

\* Retry with the same identity: beginBundle idempotent path (:137-140) then
\* Commit idempotent branch (:762-764): returns published outputs. Only re-points
\* the pointer under RepairOnIdempotent (the fix shape that does not exist today).
IdempotentRetry(e) ==
  /\ state[e] = "PUBLISHED"
  /\ RepairOnIdempotent = TRUE
  /\ pointer[e] = Nil
  /\ pointer' = [pointer EXCEPT ![e] = Present]
  /\ UNCHANGED <<state, swept>>

\* Reconcile sweep: only RUNNING/VALIDATING/FAILED (:1043); visits PUBLISHED
\* solely when SweepPublished = TRUE (counterfactual), then repairs the pointer.
Sweep(e) ==
  /\ \/ state[e] # "PUBLISHED"
     \/ SweepPublished = TRUE
  /\ swept' = [swept EXCEPT ![e] = TRUE]
  /\ \/ /\ state[e] = "PUBLISHED"
       /\ pointer' = [pointer EXCEPT ![e] = Present]
     \/ UNCHANGED pointer
  /\ UNCHANGED state

Next ==
  \/ \E e \in EXEC : Commit(e)
  \/ \E e \in EXEC : IdempotentRetry(e)
  \/ \E e \in EXEC : Sweep(e)
  \/ UNCHANGED vars

RetryProgress == \E e \in EXEC : IdempotentRetry(e)
Progress == RetryProgress \/ \E e \in EXEC : Sweep(e)

Spec ==
  /\ Init
  /\ [][Next]_vars
  /\ WF_vars(Next)
  /\ WF_vars(RetryProgress)
  /\ WF_vars(Progress)

\* Every execution that ever became PUBLISHED is eventually advertised by the
\* pointer: the two "current" surfaces (bulk-load pointer gate vs pointer-blind
\* name-latest selector) converge instead of disagreeing forever.
Agreement ==
  [](\A e \in EXEC : (state[e] = "PUBLISHED") => <>(pointer[e] = Present))

====
